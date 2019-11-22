package main

import (
	"os"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	core "k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/informers"

	// for auth with gcp
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

var (
	domainSuffix string
	kubeconfig   string
	ingressName  = "dynamic-services-ingress"
)

type listBackends struct {
	Backend []extensions.IngressBackend
}

func main() {
	//init log to JSON format
	log.SetFormatter(&log.JSONFormatter{})

	app := cli.NewApp()
	app.Name = "dynamic-ingress"
	app.EnableBashCompletion = true
	app.Usage = ""
	app.Version = "0.0.1"
	app.HelpName = "dynamic-ingress"
	app.UsageText = "dynamic-ingress [arguments...]"

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "kubeconf, c",
			Usage:       "Path to kubeconfig file",
			EnvVar:      "DYN_KUBECONFIG_PATH",
			Destination: &kubeconfig,
		},
		cli.StringFlag{
			Name:        "domain, d",
			Usage:       "Domain suffix for ingress ex: 123.example.com",
			Value:       "<namespace>.local",
			EnvVar:      "DYN_DOMAIN_SUFFIX",
			Destination: &domainSuffix,
		},
	}

	app.Action = run

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func run(c *cli.Context) error {
	var conf *rest.Config
	var err error

	if kubeconfig != "" {
		conf, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		conf, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Fatal(err.Error())
		return err
	}

	client, err := kubernetes.NewForConfig(conf)
	if err != nil {
		log.Fatal(err.Error())
		return err
	}

	// tmp var for storing service to ingress
	existIngress, err := getIngress(client)
	if err != nil {
		log.Fatal(err.Error())
		return err
	}

	log.Info("Initialized ingress synced with services: ", existIngress)

	// Watch Services Event
	// TODO: Manipulate Map for each Events and update it accordingly instead of reSync every Event occur
	watchlistSvc := cache.NewListWatchFromClient(client.CoreV1().RESTClient(), "services", "",
		fields.Everything())
	_, controllerSvc := cache.NewInformer(
		watchlistSvc,
		&core.Service{},
		time.Second*0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				svc := obj.(*core.Service)
				log.Info("Service added: ", svc.Name, " on namespace: ", svc.Namespace)
				lb := svc.Labels
				if val, found := lb["dynamic-ingress/auto"]; found {
					if val == "enabled" {
						syncIngressSvc(client)
					}
				}
			},
			DeleteFunc: func(obj interface{}) {
				svc := obj.(*core.Service)
				log.Info("Service deleted: ", svc.Name, " on namespace: ", svc.Namespace)
				lb := svc.Labels
				if _, found := lb["dynamic-ingress/auto"]; found {
					syncIngressSvc(client)
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldSvc := oldObj.(*core.Service)
				newSvc := newObj.(*core.Service)
				log.Info("Service changed: ", newSvc.Name, " on namespace: ", newSvc.Namespace)
				if _, found := oldSvc.Labels["dynamic-ingress/auto"]; found {
					syncIngressSvc(client)
				}
				if _, found := newSvc.Labels["dynamic-ingress/auto"]; found {
					syncIngressSvc(client)
				}
			},
		},
	)


	//Watch Namespace Events
	factory := informers.NewSharedInformerFactory(client, 0)
    informer := factory.Core().V1().Namespaces().Informer()
    informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc: func(obj interface{}) {
			nms := obj.(*core.Namespace)
			log.Info("Namespace added: ", nms.Name)
			lb := nms.Labels
			if _, found := lb["dynamic-ingress/auto"]; found {
					syncIngressSvc(client)
			}
		},
		DeleteFunc: func(obj interface{}) {
			nms := obj.(*core.Namespace)
			log.Info("Namespace deleted: ", nms.Name)
			lb := nms.Labels
			if _, found := lb["dynamic-ingress/auto"]; found {
				syncIngressSvc(client)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNms := oldObj.(*core.Namespace)
			newNms := newObj.(*core.Namespace)
			log.Info("Namespace changed: ", newNms.Name)
			if _, found := oldNms.Labels["dynamic-ingress/auto"]; found {
				syncIngressSvc(client)
			}
			if _, found := newNms.Labels["dynamic-ingress/auto"]; found {
				syncIngressSvc(client)
			}
		},
	})

	stopSvc := make(chan struct{})
	stopNms := make(chan struct{})
    go informer.Run(stopNms)
    if !cache.WaitForCacheSync(stopNms, informer.HasSynced) {
        log.Error("Timed out waiting for caches to sync")
    }
	go controllerSvc.Run(stopSvc)
	for {
		time.Sleep(time.Second)
	}

	return nil
}

func getNamespaceWithLabels(clientset *kubernetes.Clientset) []core.Namespace {
	var res []core.Namespace
	namespaces, err := clientset.CoreV1().Namespaces().List(metav1.ListOptions{})
	if err != nil {
		log.Error(err.Error())
		return nil
	}

	_ = len(namespaces.Items)
	for _, n := range namespaces.Items {
		lb := n.GetLabels()
		if val, inFound := lb["dynamic-ingress/auto"]; inFound {
			if val == "enabled" {
				res = append(res, n)
			}
		}
	}

	return res
}

func getSvcIngress(clientset *kubernetes.Clientset) ([]extensions.Ingress, error) {
	var resIngress []extensions.Ingress
	var listIngress map[string]listBackends
	listIngress = make(map[string]listBackends)

	// tmp get namespaces labels
	enabledNamespaces := getNamespaceWithLabels(clientset)

	log.Info("enabled NS: ", enabledNamespaces)

	// get services from all namespaces
	svc, err := clientset.CoreV1().Services("").List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// lookup from services and catch if have label "dynamic-ingress/auto: enabled" but haven't added to the ingress yet
	_ = len(svc.Items)
	for _, s := range svc.Items {
		lb := s.GetLabels()
		svcName := s.GetName()
		namespace := s.Namespace
		var backends listBackends = listIngress[namespace]
		var port int32 = 80
		if len(s.Spec.Ports) > 0 {
			port = int32(s.Spec.Ports[0].Port)
		} else {
			log.Error("Port not found for service", svcName)
			continue
		}
		if found := findB(listIngress[namespace].Backend, svcName); !found {
			add := false
			val, inFound := lb["dynamic-ingress/auto"]
			val2, inFound2 := findN(enabledNamespaces, namespace)
			if (inFound2 && val2 == "enabled") {
				if inFound && val == "disabled" {
					add = false
				}else{
					add = true
				}
			}
			if inFound && val == "enabled" {
				add = true
			}

			log.Info("svc: ", svcName, val)

			if add {
				newSvc := extensions.IngressBackend{
					ServiceName: svcName,
					ServicePort: intstr.FromInt(int(port)),
				}
				backends.Backend = append(backends.Backend, newSvc)
				listIngress[namespace] = backends
			}
		}
	}
	resIngress, err = buildIngress(listIngress)

	return resIngress, nil
}

func getIngress(clientset *kubernetes.Clientset) ([]extensions.Ingress, error) {
	var resIngress []extensions.Ingress
	var listIngress map[string]listBackends
	listIngress = make(map[string]listBackends)

	//get ingresses from all namespaces
	ing, err := clientset.ExtensionsV1beta1().Ingresses("").List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	// list all service that already on ingress
	_ = len(ing.Items)
	for _, i := range ing.Items {
		namespace := i.Namespace
		rules := i.Spec.Rules
		var backends listBackends = listIngress[namespace]
		_ = len(rules)
		for _, r := range rules {
			paths := r.HTTP.Paths
			_ = len(paths)
			for _, s := range paths {
				svcName := s.Backend.ServiceName
				if found := findB(listIngress[namespace].Backend, svcName); !found {
					backends.Backend = append(backends.Backend, s.Backend)
					listIngress[namespace] = backends
				}
			}
		}
	}
	resIngress, err = buildIngress(listIngress)

	return resIngress, nil
}

func syncIngressSvc(clientset *kubernetes.Clientset) {
	log.Info("Syncing Ingress with Service...")
	// tmp var for storing service to ingress
	svcIngr, err := getSvcIngress(clientset)
	if err != nil {
		log.Fatal(err.Error())
	}

	// tmp var for storing existing ingress
	existIngr, err := getIngress(clientset)
	if err != nil {
		log.Fatal(err.Error())
	}

	//Cleanup unused Ingress
	cleanupIngress(clientset, existIngr, svcIngr)

	// Upsert ingress to Kubernetes API
	upsertIngress(clientset, svcIngr)
}

func cleanupIngress(clientset *kubernetes.Clientset, existIngress []extensions.Ingress, svcIngress []extensions.Ingress) {
	var unusedIngress []extensions.Ingress
	_ = len(existIngress)
	for _, val := range existIngress {
		if found := findI(svcIngress, val.Name, val.Namespace); !found {
			log.Info("unusedIngress: ", unusedIngress)
			unusedIngress = append(unusedIngress, val)
		}
	}

	_ = len (unusedIngress)
	for _, val := range unusedIngress {
		log.Info("This unused ingress %s on namespace %s will be deleted", val.Name, val.Namespace)
		err := clientset.ExtensionsV1beta1().Ingresses(val.Namespace).Delete(val.Name, &metav1.DeleteOptions{})
		if err != nil {
			log.Error(err.Error())
		}
	}

}
func upsertIngress(clientset *kubernetes.Clientset, ings []extensions.Ingress) {
	_ = len(ings)
	for _, val := range ings {
		_, err := clientset.ExtensionsV1beta1().Ingresses(val.Namespace).Update(&val)
		if err != nil {
			if k8sErrors.IsNotFound(err) {
				_, err := clientset.ExtensionsV1beta1().Ingresses(val.Namespace).Create(&val)
				if err != nil {
					log.Error(err.Error())
				} else {
					log.Info("Ingress: ", val.Name, " on namespace ", val.Namespace, " created.")
				}
			} else {
				log.Error(err.Error())
			}
		} else {
			log.Info("Ingress: ", val.Name, " on namespace ", val.Namespace, " updated.")
		}
	}
}

func buildIngress(ing map[string]listBackends) ([]extensions.Ingress, error) {
	var listIngress []extensions.Ingress
	var annot map[string]string
	annot = make(map[string]string)
	annot["kubernetes.io/ingress.class"] = "haproxy"

	_ = len(ing)
	for namespace, backend := range ing {
		_ = len(backend.Backend)
		var rules []extensions.IngressRule
		for _, svc := range backend.Backend {
			domainName := svc.ServiceName + "." + genDomain(namespace)
			newRule := extensions.IngressRule{
				Host: domainName,
				IngressRuleValue: extensions.IngressRuleValue{
					HTTP: &extensions.HTTPIngressRuleValue{
						Paths: []extensions.HTTPIngressPath{
							{
								Path:    "/",
								Backend: svc,
							},
						},
					},
				},
			}
			rules = append(rules, newRule)
		}
		ingress := extensions.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:        ingressName,
				Namespace:   namespace,
				Annotations: annot,
			},
			Spec: extensions.IngressSpec{
				Rules: rules,
			},
		}
		listIngress = append(listIngress, ingress)
	}

	return listIngress, nil
}
